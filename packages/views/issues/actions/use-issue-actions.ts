"use client";

import { useCallback } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import type { Issue, UpdateIssueRequest } from "@multica/core/types";
import { api } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { useModalStore } from "@multica/core/modals";
import { useUpdateIssue } from "@multica/core/issues/mutations";
import { issueKeys } from "@multica/core/issues/queries";
import { useCreatePlan } from "@multica/core/plans/mutations";
import { agentListOptions } from "@multica/core/workspace/queries";
import { pinListOptions, useCreatePin, useDeletePin } from "@multica/core/pins";
import { useNavigation } from "../../navigation";
import { useT } from "../../i18n";

const BACKLOG_HINT_LS_KEY = "multica:backlog-agent-hint-dismissed";
const INTERNAL_PLANNER_AGENT_NAME = "规划Agent";

export interface UseIssueActionsResult {
  isPinned: boolean;
  updateField: (updates: Partial<UpdateIssueRequest>) => void;
  rerunIssue: () => void;
  togglePin: () => void;
  copyLink: () => Promise<void>;
  openCreateSubIssue: () => void;
  openSetParent: () => void;
  openAddChild: () => void;
  openDeleteConfirm: (opts?: { onDeletedNavigateTo?: string }) => void;
}

/**
 * Accepts a nullable issue so callers can invoke the hook before they've
 * early-returned on a missing issue. Returned handlers are safe no-ops when
 * `issue` is null.
 */
export function useIssueActions(issue: Issue | null): UseIssueActionsResult {
  const { t } = useT("issues");
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const navigation = useNavigation();
  const queryClient = useQueryClient();
  const user = useAuthStore((s) => s.user);
  const userId = user?.id;

  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { data: pinnedItems = [] } = useQuery({
    ...pinListOptions(wsId, userId ?? ""),
    enabled: !!userId,
  });

  const isPinned =
    !!issue &&
    pinnedItems.some(
      (p) => p.item_type === "issue" && p.item_id === issue.id,
    );

  const updateIssue = useUpdateIssue();
  const createPlan = useCreatePlan(wsId);
  const createPin = useCreatePin();
  const deletePin = useDeletePin();
  const openModal = useModalStore((s) => s.open);
  const rerunIssueMutation = useMutation({
    mutationFn: (id: string) => api.rerunIssue(id),
    onSuccess: (_task, id) => {
      toast.success(t(($) => $.actions.rerun_issue_enqueued));
      void queryClient.invalidateQueries({ queryKey: issueKeys.detail(wsId, id) });
      void queryClient.invalidateQueries({ queryKey: issueKeys.tasks(id) });
      void queryClient.invalidateQueries({ queryKey: issueKeys.tasksAll() });
      void queryClient.invalidateQueries({ queryKey: issueKeys.all(wsId) });
    },
    onError: (error) => {
      toast.error(
        error instanceof Error
          ? error.message
          : t(($) => $.actions.rerun_issue_failed),
      );
    },
  });

  const issueId = issue?.id ?? null;
  const issueTitle = issue?.title ?? "";
  const issueDescription = issue?.description ?? null;
  const issueStatus = issue?.status ?? null;
  const issueIdentifier = issue?.identifier ?? null;
  const issueProjectId = issue?.project_id ?? null;

  const updateField = useCallback(
    (updates: Partial<UpdateIssueRequest>) => {
      if (!issueId) return;
      const plannerAgent =
        updates.assignee_type === "agent" && updates.assignee_id
          ? agents.find(
              (agent) =>
                agent.id === updates.assignee_id &&
                !agent.archived_at &&
                agent.is_internal &&
                agent.name === INTERNAL_PLANNER_AGENT_NAME,
            )
          : undefined;
      if (plannerAgent) {
        const prompt = [issueTitle, issueDescription]
          .map((part) => part?.trim() ?? "")
          .filter(Boolean)
          .join("\n\n");
        createPlan.mutate(
          {
            title: issueTitle.trim() || undefined,
            prompt,
            planner_agent_id: plannerAgent.id,
            project_id: issueProjectId,
            source_issue_id: issueId,
          },
          {
            onSuccess: (plan) => {
              toast.success(t(($) => $.detail.plan_created), {
                action: {
                  label: t(($) => $.detail.open_plan),
                  onClick: () => navigation.push(paths.planDetail(plan.id)),
                },
              });
            },
            onError: (error) =>
              toast.error(
                error instanceof Error
                  ? error.message
                  : t(($) => $.detail.plan_create_failed),
              ),
          },
        );
        return;
      }
      updateIssue.mutate(
        { id: issueId, ...updates },
        {
          onError: (err) =>
            toast.error(
              err instanceof Error && err.message
                ? err.message
                : t(($) => $.detail.update_failed),
            ),
        },
      );
      // Hint: assigning an agent to a backlog issue won't trigger execution
      // until the issue is moved to an active status.
      if (
        updates.assignee_type === "agent" &&
        updates.assignee_id &&
        issueStatus === "backlog" &&
        typeof window !== "undefined" &&
        localStorage.getItem(BACKLOG_HINT_LS_KEY) !== "true"
      ) {
        openModal("issue-backlog-agent-hint", { issueId });
      }
    },
    [
      issueId,
      issueTitle,
      issueDescription,
      issueStatus,
      issueProjectId,
      agents,
      createPlan,
      updateIssue,
      openModal,
      navigation,
      paths,
      t,
    ],
  );

  const togglePin = useCallback(() => {
    if (!issueId) return;
    if (isPinned) {
      deletePin.mutate({ itemType: "issue", itemId: issueId });
    } else {
      createPin.mutate({ item_type: "issue", item_id: issueId });
    }
  }, [isPinned, issueId, createPin, deletePin]);

  const rerunIssue = useCallback(() => {
    if (!issueId) return;
    rerunIssueMutation.mutate(issueId);
  }, [issueId, rerunIssueMutation]);

  const copyLink = useCallback(async () => {
    if (!issueId) return;
    const url = navigation.getShareableUrl(paths.issueDetail(issueId));
    try {
      await navigator.clipboard.writeText(url);
      toast.success(t(($) => $.detail.link_copied));
    } catch {
      toast.error(t(($) => $.detail.link_copy_failed));
    }
  }, [paths, issueId, navigation, t]);

  const openCreateSubIssue = useCallback(() => {
    if (!issueId) return;
    openModal("create-issue", {
      parent_issue_id: issueId,
      parent_issue_identifier: issueIdentifier,
      ...(issueProjectId ? { project_id: issueProjectId } : {}),
    });
  }, [openModal, issueId, issueIdentifier, issueProjectId]);

  const openSetParent = useCallback(() => {
    if (!issueId) return;
    openModal("issue-set-parent", { issueId });
  }, [openModal, issueId]);

  const openAddChild = useCallback(() => {
    if (!issueId) return;
    openModal("issue-add-child", { issueId });
  }, [openModal, issueId]);

  const openDeleteConfirm = useCallback(
    (opts?: { onDeletedNavigateTo?: string }) => {
      if (!issueId) return;
      openModal("issue-delete-confirm", {
        issueId,
        identifier: issueIdentifier,
        onDeletedNavigateTo: opts?.onDeletedNavigateTo,
      });
    },
    [openModal, issueId, issueIdentifier],
  );

  return {
    isPinned,
    updateField,
    rerunIssue,
    togglePin,
    copyLink,
    openCreateSubIssue,
    openSetParent,
    openAddChild,
    openDeleteConfirm,
  };
}
