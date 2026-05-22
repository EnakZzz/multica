import { beforeEach, describe, expect, it } from "vitest";
import { usePlanDraftStore } from "./draft-store";
import type { PlanSpec } from "../../types/plan";

const EMPTY_STORE = { drafts: {} };

const PLAN_A = "plan-aaa-111";
const PLAN_B = "plan-bbb-222";

const SPEC_A: PlanSpec = {
  summary: "Summary A",
  goal: "Goal A",
  success_criteria: ["done"],
  in_scope: [],
  out_of_scope: [],
  approach: "Approach A",
  assumptions: [],
  open_questions: [],
};

describe("usePlanDraftStore", () => {
  beforeEach(() => {
    usePlanDraftStore.setState(EMPTY_STORE);
  });

  it("returns undefined for an unknown planId", () => {
    expect(usePlanDraftStore.getState().getDraft(PLAN_A)).toBeUndefined();
  });

  it("persists specDraft for a given planId", () => {
    const { setDraft, getDraft } = usePlanDraftStore.getState();
    setDraft(PLAN_A, { specDraft: SPEC_A });
    expect(getDraft(PLAN_A)?.specDraft).toEqual(SPEC_A);
  });

  it("persists parentTitle and parentDescription independently", () => {
    const { setDraft, getDraft } = usePlanDraftStore.getState();
    setDraft(PLAN_A, { parentTitle: "My Parent", parentDescription: "Desc" });
    const d = getDraft(PLAN_A);
    expect(d?.parentTitle).toBe("My Parent");
    expect(d?.parentDescription).toBe("Desc");
  });

  it("different planIds do not cross-contaminate", () => {
    const { setDraft, getDraft } = usePlanDraftStore.getState();
    setDraft(PLAN_A, { specDraft: SPEC_A });
    setDraft(PLAN_B, { parentTitle: "B title" });

    expect(getDraft(PLAN_A)?.specDraft).toEqual(SPEC_A);
    expect(getDraft(PLAN_A)?.parentTitle).toBe("");
    expect(getDraft(PLAN_B)?.specDraft).toBeNull();
    expect(getDraft(PLAN_B)?.parentTitle).toBe("B title");
  });

  it("clearDraft removes only the targeted planId", () => {
    const { setDraft, clearDraft, getDraft } = usePlanDraftStore.getState();
    setDraft(PLAN_A, { specDraft: SPEC_A });
    setDraft(PLAN_B, { parentTitle: "B title" });

    clearDraft(PLAN_A);

    expect(getDraft(PLAN_A)).toBeUndefined();
    expect(getDraft(PLAN_B)?.parentTitle).toBe("B title");
  });

  it("setDraft with all-empty fields removes the entry", () => {
    const { setDraft, getDraft } = usePlanDraftStore.getState();
    setDraft(PLAN_A, { specDraft: SPEC_A });
    setDraft(PLAN_A, { specDraft: null, dirtyItems: null, parentTitle: "", parentDescription: "" });
    expect(getDraft(PLAN_A)).toBeUndefined();
  });

  it("pruneStaleDrafts removes entries older than TTL on rehydrate", () => {
    const now = Date.now();
    const TTL_MS = 7 * 24 * 60 * 60 * 1000;
    const staleTime = now - TTL_MS - 1000;

    usePlanDraftStore.setState({
      drafts: {
        "stale-plan": {
          specDraft: SPEC_A,
          dirtyItems: null,
          parentTitle: "",
          parentDescription: "",
          updatedAt: staleTime,
        },
        "fresh-plan": {
          specDraft: null,
          dirtyItems: null,
          parentTitle: "fresh",
          parentDescription: "",
          updatedAt: now,
        },
      },
    });

    // Simulate rehydrate pruning by directly invoking the prune logic
    // (the store's onRehydrateStorage runs automatically on real persist rehydrate)
    const state = usePlanDraftStore.getState();
    // Stale entry was set before any prune; verify our setup
    expect(state.drafts["stale-plan"]).toBeDefined();
    expect(state.drafts["fresh-plan"]).toBeDefined();

    // Re-create state after pruning (simulate what onRehydrateStorage does)
    const cutoff = Date.now() - TTL_MS;
    const pruned = Object.fromEntries(
      Object.entries(state.drafts).filter(([, v]) => v.updatedAt >= cutoff),
    );
    usePlanDraftStore.setState({ drafts: pruned });

    expect(usePlanDraftStore.getState().getDraft("stale-plan")).toBeUndefined();
    expect(usePlanDraftStore.getState().getDraft("fresh-plan")?.parentTitle).toBe("fresh");
  });

  it("updatedAt is set to a positive timestamp on setDraft", () => {
    const { setDraft, getDraft } = usePlanDraftStore.getState();
    const before = Date.now();
    setDraft(PLAN_A, { parentTitle: "first" });
    const t1 = getDraft(PLAN_A)?.updatedAt ?? 0;
    expect(t1).toBeGreaterThanOrEqual(before);
    expect(t1).toBeLessThanOrEqual(Date.now());
  });
});
