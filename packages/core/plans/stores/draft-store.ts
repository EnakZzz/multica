import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import { createWorkspaceAwareStorage, registerForWorkspaceRehydration } from "../../platform/workspace-storage";
import { defaultStorage } from "../../platform/storage";
import type { PlanItem, PlanSpec } from "../../types/plan";

/**
 * Per-plan draft persistence for unsaved spec edits, task item edits, and
 * parent issue fields. Survives route navigation (unmount/remount) without
 * requiring an explicit server-side "save draft" API.
 *
 * Keys are plan-scoped. createWorkspaceAwareStorage already partitions by
 * workspace slug, so a planId collision across workspaces is impossible in
 * practice — but the workspace scope gives an extra safety net.
 *
 * Clear triggers (product expectation):
 *   - Successful save (updatePlan / approvePlanSpec)
 *   - Successful commit (commitPlan)
 *   - Caller explicitly calls clearDraft(planId)
 *
 * Drafts older than 7 days are pruned on store init to prevent unbounded growth.
 */

export interface PlanDraft {
  specDraft: PlanSpec | null;
  dirtyItems: PlanItem[] | null;
  parentTitle: string;
  parentDescription: string;
  updatedAt: number;
}

interface PlanDraftStore {
  drafts: Record<string, PlanDraft>;
  getDraft: (planId: string) => PlanDraft | undefined;
  setDraft: (planId: string, patch: Partial<Omit<PlanDraft, "updatedAt">>) => void;
  clearDraft: (planId: string) => void;
}

const TTL_MS = 7 * 24 * 60 * 60 * 1000;

function pruneStaleDrafts(drafts: Record<string, PlanDraft>): Record<string, PlanDraft> {
  const cutoff = Date.now() - TTL_MS;
  const out: Record<string, PlanDraft> = {};
  for (const [k, v] of Object.entries(drafts)) {
    if (v.updatedAt >= cutoff) {
      out[k] = v;
    }
  }
  return out;
}

function isEmptyDraft(draft: PlanDraft): boolean {
  return (
    draft.specDraft === null &&
    draft.dirtyItems === null &&
    draft.parentTitle === "" &&
    draft.parentDescription === ""
  );
}

export const usePlanDraftStore = create<PlanDraftStore>()(
  persist(
    (set, get) => ({
      drafts: {},
      getDraft: (planId) => get().drafts[planId],
      setDraft: (planId, patch) =>
        set((s) => {
          const existing = s.drafts[planId] ?? {
            specDraft: null,
            dirtyItems: null,
            parentTitle: "",
            parentDescription: "",
            updatedAt: 0,
          };
          const next: PlanDraft = { ...existing, ...patch, updatedAt: Date.now() };
          if (isEmptyDraft(next)) {
            // Don't persist empty drafts
            if (!(planId in s.drafts)) return s;
            const nextDrafts = { ...s.drafts };
            delete nextDrafts[planId];
            return { drafts: nextDrafts };
          }
          return { drafts: { ...s.drafts, [planId]: next } };
        }),
      clearDraft: (planId) =>
        set((s) => {
          if (!(planId in s.drafts)) return s;
          const next = { ...s.drafts };
          delete next[planId];
          return { drafts: next };
        }),
    }),
    {
      name: "multica_plan_drafts",
      storage: createJSONStorage(() => createWorkspaceAwareStorage(defaultStorage)),
      onRehydrateStorage: () => (state) => {
        if (state) {
          state.drafts = pruneStaleDrafts(state.drafts);
        }
      },
    },
  ),
);

registerForWorkspaceRehydration(() => usePlanDraftStore.persist.rehydrate());
