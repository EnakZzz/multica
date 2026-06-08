"use client";

import { useState } from "react";
import { CheckCircle2, FileText, Upload, X } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@multica/ui/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import { useWorkspaceId } from "@multica/core/hooks";
import {
  useImportPipelineYaml,
  useValidatePipelineYamlImport,
} from "@multica/core/pipelines/mutations";
import type {
  Pipeline,
  PipelineImportValidationResponse,
} from "@multica/core/types";

export function PipelineImportDialog({
  open,
  onOpenChange,
  pipelineId,
  onImported,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  pipelineId?: string;
  onImported?: (pipeline: Pipeline) => void;
}) {
  const wsId = useWorkspaceId();
  const validateImport = useValidatePipelineYamlImport();
  const importPipeline = useImportPipelineYaml(wsId);
  const [content, setContent] = useState("");
  const [fileName, setFileName] = useState("");
  const [validation, setValidation] = useState<PipelineImportValidationResponse | null>(null);

  const payload = {
    content,
    pipeline_id: pipelineId ?? null,
  };

  const readFile = async (file: File | null | undefined) => {
    if (!file) return;
    const name = file.name.trim();
    if (!/\.(ya?ml)$/i.test(name)) {
      toast.error("Select a .yaml or .yml file");
      return;
    }
    try {
      const text = await file.text();
      setContent(text);
      setFileName(name);
      setValidation(null);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Failed to read YAML file");
    }
  };

  const clearFile = () => {
    setContent("");
    setFileName("");
    setValidation(null);
  };

  const validate = async () => {
    try {
      const result = await validateImport.mutateAsync(payload);
      setValidation(result);
      if (result.valid) {
        toast.success("YAML validated");
      } else {
        toast.error(result.errors[0] ?? "Invalid workflow YAML");
      }
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Failed to validate YAML");
    }
  };

  const submit = async () => {
    try {
      const pipeline = await importPipeline.mutateAsync(payload);
      clearFile();
      onOpenChange(false);
      toast.success(pipelineId ? "Workflow imported" : "Workflow created");
      onImported?.(pipeline);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Failed to import workflow");
    }
  };

  const errors = validation?.valid === false ? validation.errors : [];

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-3xl">
        <DialogHeader>
          <DialogTitle>Import YAML</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <label
            className="flex min-h-48 cursor-pointer flex-col items-center justify-center gap-3 rounded-md border border-dashed border-border bg-muted/20 px-4 py-8 text-center transition-colors hover:bg-muted/40"
            onDragOver={(event) => {
              event.preventDefault();
            }}
            onDrop={(event) => {
              event.preventDefault();
              void readFile(event.dataTransfer.files.item(0));
            }}
          >
            <input
              type="file"
              accept=".yaml,.yml,text/yaml,application/x-yaml"
              aria-label="Choose YAML file"
              className="sr-only"
              onChange={(event) => {
                void readFile(event.target.files?.item(0));
                event.target.value = "";
              }}
            />
            <span className="flex h-11 w-11 items-center justify-center rounded-md border bg-background">
              <Upload className="h-5 w-5 text-muted-foreground" />
            </span>
            <span className="text-sm font-medium">Choose a YAML file</span>
            <span className="text-xs text-muted-foreground">.yaml or .yml</span>
          </label>
          {fileName && (
            <div className="flex items-center gap-2 rounded-md border bg-muted/20 px-3 py-2 text-sm">
              <FileText className="h-4 w-4 text-muted-foreground" />
              <span className="min-w-0 flex-1 truncate">{fileName}</span>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className="h-7 w-7"
                onClick={clearFile}
                title="Remove file"
              >
                <X className="h-4 w-4" />
              </Button>
            </div>
          )}
          {validation?.valid && validation.pipeline && (
            <div className="flex items-center gap-2 rounded-md border border-emerald-500/40 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-700 dark:text-emerald-300">
              <CheckCircle2 className="h-4 w-4" />
              <span className="truncate">
                {validation.pipeline.name} - {validation.pipeline.nodes.length} nodes
              </span>
            </div>
          )}
          {errors.length > 0 && (
            <div className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
              {errors[0]}
            </div>
          )}
          <div className="flex justify-end gap-2">
            <Button variant="ghost" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button
              variant="outline"
              onClick={validate}
              disabled={!content.trim() || validateImport.isPending}
            >
              Validate
            </Button>
            <Button
              onClick={submit}
              disabled={!content.trim() || importPipeline.isPending}
            >
              <Upload className="mr-1 h-4 w-4" />
              Import
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
