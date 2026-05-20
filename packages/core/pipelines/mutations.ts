import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { issueKeys } from "../issues/queries";
import type {
  CreatePipelineRequest,
  DuplicatePipelineRequest,
  ImportPipelineYamlRequest,
  RunPipelineRequest,
  UpdatePipelineRequest,
} from "../types";
import { pipelineKeys } from "./queries";

export function useCreatePipeline(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: CreatePipelineRequest) => api.createPipeline(data),
    onSuccess: (pipeline) => {
      qc.invalidateQueries({ queryKey: pipelineKeys.list(wsId) });
      qc.setQueryData(pipelineKeys.detail(wsId, pipeline.id), pipeline);
    },
  });
}

export function useUpdatePipeline(wsId: string, pipelineId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: UpdatePipelineRequest) => api.updatePipeline(pipelineId, data),
    onSuccess: (pipeline) => {
      qc.setQueryData(pipelineKeys.detail(wsId, pipeline.id), pipeline);
      qc.invalidateQueries({ queryKey: pipelineKeys.list(wsId) });
    },
  });
}

export function useDeletePipeline(wsId: string, pipelineId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.deletePipeline(pipelineId),
    onSuccess: () => {
      qc.removeQueries({ queryKey: pipelineKeys.detail(wsId, pipelineId) });
      qc.invalidateQueries({ queryKey: pipelineKeys.list(wsId) });
    },
  });
}

export function useDuplicatePipeline(wsId: string, pipelineId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data?: DuplicatePipelineRequest) => api.duplicatePipeline(pipelineId, data ?? {}),
    onSuccess: (pipeline) => {
      qc.invalidateQueries({ queryKey: pipelineKeys.list(wsId) });
      qc.setQueryData(pipelineKeys.detail(wsId, pipeline.id), pipeline);
    },
  });
}

export function useValidatePipelineYamlImport() {
  return useMutation({
    mutationFn: (data: ImportPipelineYamlRequest) => api.validatePipelineYamlImport(data),
  });
}

export function useImportPipelineYaml(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: ImportPipelineYamlRequest) => api.importPipelineYaml(data),
    onSuccess: (pipeline) => {
      qc.invalidateQueries({ queryKey: pipelineKeys.list(wsId) });
      qc.setQueryData(pipelineKeys.detail(wsId, pipeline.id), pipeline);
    },
  });
}

export function useRunPipeline(wsId: string, pipelineId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data?: RunPipelineRequest) => api.runPipeline(pipelineId, data ?? {}),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: issueKeys.all(wsId) });
      qc.invalidateQueries({ queryKey: pipelineKeys.detail(wsId, pipelineId) });
    },
  });
}
