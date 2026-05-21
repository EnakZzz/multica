import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { issueKeys } from "../issues/queries";
import type { ApprovePlanSpecRequest, ClarifyPlanSpecRequest, CommitPlanRequest, CreatePlanRequest, UpdatePlanRequest } from "../types";
import { planKeys } from "./queries";

export function useCreatePlan(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: CreatePlanRequest) => api.createPlan(data),
    onSuccess: (plan) => {
      qc.invalidateQueries({ queryKey: planKeys.list(wsId) });
      qc.setQueryData(planKeys.detail(wsId, plan.id), plan);
    },
  });
}

export function useUpdatePlan(wsId: string, planId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: UpdatePlanRequest) => api.updatePlan(planId, data),
    onSuccess: (plan) => {
      qc.setQueryData(planKeys.detail(wsId, plan.id), plan);
      qc.invalidateQueries({ queryKey: planKeys.list(wsId) });
    },
  });
}

export function useRerunPlan(wsId: string, planId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.rerunPlan(planId),
    onSuccess: (plan) => {
      qc.setQueryData(planKeys.detail(wsId, plan.id), plan);
      qc.invalidateQueries({ queryKey: planKeys.list(wsId) });
    },
  });
}

export function useApprovePlanSpec(wsId: string, planId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: ApprovePlanSpecRequest = {}) => api.approvePlanSpec(planId, data),
    onSuccess: (plan) => {
      qc.setQueryData(planKeys.detail(wsId, plan.id), plan);
      qc.invalidateQueries({ queryKey: planKeys.list(wsId) });
    },
  });
}

export function useClarifyPlanSpec(wsId: string, planId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: ClarifyPlanSpecRequest) => api.clarifyPlanSpec(planId, data),
    onSuccess: (plan) => {
      qc.setQueryData(planKeys.detail(wsId, plan.id), plan);
      qc.invalidateQueries({ queryKey: planKeys.list(wsId) });
    },
  });
}

export function useCommitPlan(wsId: string, planId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: CommitPlanRequest = {}) => api.commitPlan(planId, data),
    onSuccess: (plan) => {
      qc.setQueryData(planKeys.detail(wsId, plan.id), plan);
      qc.invalidateQueries({ queryKey: planKeys.list(wsId) });
      qc.invalidateQueries({ queryKey: issueKeys.all(wsId) });
      if (plan.parent_issue_id) {
        qc.invalidateQueries({ queryKey: issueKeys.children(wsId, plan.parent_issue_id) });
      }
    },
  });
}
