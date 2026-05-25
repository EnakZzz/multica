import { afterEach, describe, expect, it, vi } from "vitest";
import { z } from "zod";
import { ApiClient } from "./client";
import { parseWithFallback } from "./schema";

// Helper: stub fetch with a single JSON response. Status defaults to 200.
function stubFetchJson(body: unknown, status = 200) {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      new Response(typeof body === "string" ? body : JSON.stringify(body), {
        status,
        headers: { "Content-Type": "application/json" },
      }),
    ),
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
});

// These tests cover the five failure modes that white-screened the desktop
// app in past incidents. The contract is: a malformed response degrades to
// an empty/safe shape, never throws into React.
describe("ApiClient schema fallback", () => {
  describe("listTimeline", () => {
    it("falls back to an empty array when the body is null", async () => {
      stubFetchJson(null);
      const client = new ApiClient("https://api.example.test");
      const entries = await client.listTimeline("issue-1");
      expect(entries).toEqual([]);
    });

    it("falls back when the body is not an array", async () => {
      stubFetchJson({ wrong: "shape" });
      const client = new ApiClient("https://api.example.test");
      const entries = await client.listTimeline("issue-1");
      expect(entries).toEqual([]);
    });

    it("accepts a new entry type rather than crashing on enum drift", async () => {
      stubFetchJson([
        {
          type: "future_kind", // not in TS union
          id: "e-1",
          actor_type: "member",
          actor_id: "u-1",
          created_at: "2026-01-01T00:00:00Z",
        },
      ]);
      const client = new ApiClient("https://api.example.test");
      const entries = await client.listTimeline("issue-1");
      expect(entries).toHaveLength(1);
      expect(entries[0]?.type).toBe("future_kind");
    });

    // Forward-compat: when the server adds a new field to an existing
    // shape, `.loose()` lets it pass through unchanged. Without `.loose()`
    // zod 4 strips it, which would silently break a future TS type that
    // adopts the field — see schemas.ts header comment.
    it("preserves unknown fields the schema didn't list", async () => {
      stubFetchJson([
        {
          type: "comment",
          id: "e-1",
          actor_type: "member",
          actor_id: "u-1",
          created_at: "2026-01-01T00:00:00Z",
          // New server-side field not present in TimelineEntrySchema:
          future_field: { nested: "value" },
        },
      ]);
      const client = new ApiClient("https://api.example.test");
      const entries = await client.listTimeline("issue-1");
      const entry = entries[0] as unknown as Record<string, unknown>;
      expect(entry.future_field).toEqual({ nested: "value" });
    });
  });

  describe("listIssues", () => {
    it("falls back to an empty list when the response is malformed", async () => {
      // `issues` having the wrong type triggers the fallback. An object
      // with only unexpected keys would *succeed* parsing now (every
      // declared field has a default) and just pass the extras through
      // via `.loose()`, so we use a wrong-type payload here instead.
      stubFetchJson({ issues: "not-an-array", total: 0 });
      const client = new ApiClient("https://api.example.test");
      const res = await client.listIssues();
      expect(res).toEqual({ issues: [], total: 0 });
    });

    it("uses safe unit test defaults for legacy issue responses", async () => {
      stubFetchJson({
        id: "issue-1",
        workspace_id: "ws-1",
        number: 1,
        identifier: "MUL-1",
        title: "Legacy issue",
        description: null,
        status: "todo",
        priority: "none",
        assignee_type: null,
        assignee_id: null,
        creator_type: "member",
        creator_id: "user-1",
        parent_issue_id: null,
        project_id: null,
        position: 0,
        start_date: null,
        due_date: null,
        created_at: "2026-05-21T00:00:00Z",
        updated_at: "2026-05-21T00:00:00Z",
      });
      const client = new ApiClient("https://api.example.test");
      const issue = await client.getIssue("issue-1");
      expect(issue.unit_test_checklist).toEqual([]);
      expect(issue.unit_test_status).toBe("not_required");
      expect(issue.unit_test_iteration_count).toBe(0);
      expect(issue.unit_test_max_iterations).toBe(2);
    });

    it("parses issue unit test checklist result fields", async () => {
      stubFetchJson({
        id: "issue-1",
        workspace_id: "ws-1",
        number: 1,
        identifier: "MUL-1",
        title: "Checklist issue",
        description: null,
        status: "todo",
        priority: "none",
        assignee_type: "agent",
        assignee_id: "agent-1",
        creator_type: "member",
        creator_id: "user-1",
        parent_issue_id: null,
        project_id: null,
        position: 0,
        start_date: null,
        due_date: null,
        unit_test_status: "failed",
        unit_test_iteration_count: 1,
        unit_test_max_iterations: 2,
        unit_test_checklist: [
          {
            id: "check-1",
            title: "Focused unit test",
            command: "go test ./internal/service -run TestFocused -count=1",
            expected: "passes",
            required: true,
            status: "failed",
            last_run_at: "2026-05-21T00:00:00Z",
            output_excerpt: "expected pass",
            failure_summary: "test failed",
            task_id: "task-1",
          },
        ],
        created_at: "2026-05-21T00:00:00Z",
        updated_at: "2026-05-21T00:00:00Z",
      });
      const client = new ApiClient("https://api.example.test");
      const issue = await client.getIssue("issue-1");
      expect(issue.unit_test_status).toBe("failed");
      expect(issue.unit_test_checklist?.[0]).toMatchObject({
        id: "check-1",
        status: "failed",
        failure_summary: "test failed",
      });
    });
  });

  describe("listGroupedIssues", () => {
    it("falls back to empty groups when the response is malformed", async () => {
      stubFetchJson({ groups: "not-an-array" });
      const client = new ApiClient("https://api.example.test");
      const res = await client.listGroupedIssues({ group_by: "assignee" });
      expect(res).toEqual({ groups: [] });
    });
  });

  describe("quickCreateIssue", () => {
    it("accepts a plan response from planner-routed quick create", async () => {
      stubFetchJson({ plan_id: "plan-1" });
      const client = new ApiClient("https://api.example.test");
      const res = await client.quickCreateIssue({ agent_id: "agent-1", prompt: "Plan this" });
      expect(res).toEqual({ task_id: "", plan_id: "plan-1" });
    });

    it("uses safe defaults when quick create response fields are missing or malformed", async () => {
      stubFetchJson({ task_id: 123, extra: "kept" });
      const client = new ApiClient("https://api.example.test");
      const res = await client.quickCreateIssue({ agent_id: "agent-1", prompt: "Create this" });
      expect(res).toMatchObject({ task_id: "", plan_id: "" });
    });
  });

  describe("listPlans", () => {
    it("falls back to an empty plan list when the response is malformed", async () => {
      stubFetchJson({ plans: "not-an-array" });
      const client = new ApiClient("https://api.example.test");
      const res = await client.listPlans();
      expect(res).toEqual({ plans: [] });
    });

    it("uses safe spec defaults and accepts spec_review status", async () => {
      stubFetchJson({
        plans: [
          {
            id: "plan-1",
            workspace_id: "ws-1",
            title: "Plan",
            prompt: "Build it",
            status: "spec_review",
            planner_agent_id: "agent-1",
            spec: { goal: "Ship it" },
          },
        ],
      });
      const client = new ApiClient("https://api.example.test");
      const res = await client.listPlans();
      expect(res.plans[0]?.status).toBe("spec_review");
      expect(res.plans[0]?.spec).toMatchObject({
        summary: "",
        goal: "Ship it",
        success_criteria: [],
        acceptance_scenarios: [],
        design_decisions: [],
        verification_commands: [],
        open_questions: [],
        clarifications: [],
      });
      expect(res.plans[0]?.spec_approved_at).toBeNull();
      expect(res.plans[0]?.committed_spec).toBeNull();
    });

    it("parses plan spec clarification history", async () => {
      stubFetchJson({
        id: "plan-1",
        workspace_id: "ws-1",
        title: "Plan",
        prompt: "Build it",
        status: "spec_review",
        planner_agent_id: "agent-1",
        spec: {
          summary: "Draft",
          goal: "Ship it",
          acceptance_scenarios: [
            { name: "Approve", given: "Draft exists", when: "User approves", then: "Items are generated" },
          ],
          design_decisions: ["Keep approval before execution"],
          verification_commands: ["go test ./internal/handler -run TestPlan"],
          clarifications: [{ question: "Which repo?", answer: "multica" }],
        },
        committed_spec: {
          summary: "Committed draft",
          goal: "Ship committed scope",
        },
      });
      const client = new ApiClient("https://api.example.test");
      const res = await client.getPlan("plan-1");
      expect(res.spec.clarifications).toEqual([{ question: "Which repo?", answer: "multica" }]);
      expect(res.spec.acceptance_scenarios).toEqual([
        { name: "Approve", given: "Draft exists", when: "User approves", then: "Items are generated" },
      ]);
      expect(res.spec.design_decisions).toEqual(["Keep approval before execution"]);
      expect(res.spec.verification_commands).toEqual(["go test ./internal/handler -run TestPlan"]);
      expect(res.committed_spec?.summary).toBe("Committed draft");
      expect(res.committed_spec?.acceptance_scenarios).toEqual([]);
    });

    it("uses safe execution contract defaults for malformed plan items", async () => {
      stubFetchJson({
        plans: [
          {
            id: "plan-1",
            workspace_id: "ws-1",
            title: "Plan",
            prompt: "Build it",
            status: "ready",
            planner_agent_id: "agent-1",
            items: [
              {
                id: "item-1",
                plan_id: "plan-1",
                position: 1,
                title: "Build backend",
                acceptance_criteria: "not-an-array",
                suggested_test_commands: null,
                context_resources: ["server/internal/handler/plan.go"],
                risk_notes: [123],
                execution_kind: "not-valid",
                confirmation_question: null,
                confirmation_reason: 123,
                required_evidence: "not-an-array",
                depends_on_positions: "not-an-array",
              },
            ],
          },
        ],
      });
      const client = new ApiClient("https://api.example.test");
      const res = await client.listPlans();
      expect(res.plans[0]?.items[0]).toMatchObject({
        acceptance_criteria: [],
        suggested_test_commands: [],
        unit_test_checklist: [],
        context_resources: ["server/internal/handler/plan.go"],
        risk_notes: [],
        execution_kind: "agent_task",
        confirmation_question: "",
        confirmation_reason: "",
        required_evidence: [],
        requires_git_commit: true,
        branch_name: "",
        depends_on_positions: [],
      });
    });
  });

  describe("listPipelines", () => {
    it("uses schema defaults for optional pipeline fields", async () => {
      stubFetchJson({
        pipelines: [
          {
            id: "pipe-1",
            workspace_id: "ws-1",
            name: "Production",
          },
        ],
      });
      const client = new ApiClient("https://api.example.test");
      const res = await client.listPipelines();
      expect(res.total).toBe(0);
      expect(res.pipelines[0]).toMatchObject({
        id: "pipe-1",
        description: "",
        is_system: false,
        system_key: null,
        editable: true,
        deletable: true,
        nodes: [],
      });
    });

    it("parses built-in pipeline metadata safely", async () => {
      stubFetchJson({
        pipelines: [
          {
            id: "pipe-1",
            workspace_id: "ws-1",
            name: "Systematic Debugging",
            is_system: true,
            system_key: "systematic-debugging",
            editable: false,
            deletable: false,
          },
        ],
      });
      const client = new ApiClient("https://api.example.test");
      const res = await client.listPipelines();
      expect(res.pipelines[0]).toMatchObject({
        is_system: true,
        system_key: "systematic-debugging",
        editable: false,
        deletable: false,
      });
    });

    it("falls back to an empty pipeline list when the response is malformed", async () => {
      stubFetchJson({ pipelines: "not-an-array", total: 1 });
      const client = new ApiClient("https://api.example.test");
      const res = await client.listPipelines();
      expect(res).toEqual({ pipelines: [], total: 0 });
    });

    it("accepts review gate node types and downgrades unknown node types", async () => {
      stubFetchJson({
        pipelines: [
          {
            id: "pipe-1",
            workspace_id: "ws-1",
            name: "Review gates",
            nodes: [
              { id: "node-1", pipeline_id: "pipe-1", key: "spec", type: "spec_review", title: "Spec review" },
              { id: "node-2", pipeline_id: "pipe-1", key: "code", type: "code_review", title: "Code review" },
              { id: "node-3", pipeline_id: "pipe-1", key: "future", type: "future_gate", title: "Future gate" },
            ],
          },
        ],
      });
      const client = new ApiClient("https://api.example.test");
      const res = await client.listPipelines();
      expect(res.pipelines[0]?.nodes.map((node) => node.type)).toEqual(["spec_review", "code_review", "issue"]);
    });
  });

  describe("validatePipelineYamlImport", () => {
    it("uses schema defaults for missing import validation fields", async () => {
      stubFetchJson({ valid: true });
      const client = new ApiClient("https://api.example.test");
      const res = await client.validatePipelineYamlImport({ content: "name: Test" });
      expect(res).toEqual({
        valid: true,
        errors: [],
        pipeline: null,
      });
    });

    it("falls back when import validation response is malformed", async () => {
      stubFetchJson({ valid: "yes", errors: [] });
      const client = new ApiClient("https://api.example.test");
      const res = await client.validatePipelineYamlImport({ content: "name: Test" });
      expect(res).toEqual({
        valid: false,
        errors: [],
        pipeline: null,
      });
    });
  });

  describe("listComments", () => {
    it("returns [] when the response is not an array", async () => {
      stubFetchJson({ wrong: "shape" });
      const client = new ApiClient("https://api.example.test");
      const comments = await client.listComments("issue-1");
      expect(comments).toEqual([]);
    });
  });

  describe("listIssueSubscribers", () => {
    it("returns [] when the response is null", async () => {
      stubFetchJson(null);
      const client = new ApiClient("https://api.example.test");
      const subs = await client.listIssueSubscribers("issue-1");
      expect(subs).toEqual([]);
    });
  });

  describe("listChildIssues", () => {
    it("returns { issues: [] } when the issues field is missing", async () => {
      stubFetchJson({});
      const client = new ApiClient("https://api.example.test");
      const res = await client.listChildIssues("issue-1");
      expect(res).toEqual({ issues: [] });
    });
  });

  describe("listIssueDependencies", () => {
    it("falls back to empty dependency lists when the response is malformed", async () => {
      stubFetchJson({ blocked_by: "not-an-array", blocks: [] });
      const client = new ApiClient("https://api.example.test");
      const res = await client.listIssueDependencies("issue-1");
      expect(res).toEqual({ blocked_by: [], blocks: [] });
    });
  });

  // Agent template catalog is hit by the desktop create-agent picker.
  // Installed desktop builds outlive any given server, so the shape MUST
  // survive future field renames / wrapping without crashing. Each test
  // here mirrors a concrete future drift we want to absorb.
  describe("listAgentTemplates", () => {
    it("falls back to [] when the body is null", async () => {
      stubFetchJson(null);
      const client = new ApiClient("https://api.example.test");
      const tmpls = await client.listAgentTemplates();
      expect(tmpls).toEqual([]);
    });

    it("defaults skills to [] when the field is missing from a template", async () => {
      // Future server: drops `skills` because the picker no longer reads
      // them. Picker code calls `template.skills.length` — must not throw.
      stubFetchJson([{ slug: "x", name: "X" }]);
      const client = new ApiClient("https://api.example.test");
      const tmpls = await client.listAgentTemplates();
      expect(tmpls).toHaveLength(1);
      expect(tmpls[0]?.skills).toEqual([]);
    });

    it("accepts the bare-array shape (current contract)", async () => {
      stubFetchJson([
        { slug: "a", name: "A", description: "", skills: [] },
        { slug: "b", name: "B", description: "", skills: [] },
      ]);
      const client = new ApiClient("https://api.example.test");
      const tmpls = await client.listAgentTemplates();
      expect(tmpls.map((t) => t.slug)).toEqual(["a", "b"]);
    });

    it("accepts a future {templates: [...]} envelope without breaking", async () => {
      // Server migrates to a paginated envelope. We unwrap so the picker
      // keeps working on the older bare-array consumer.
      stubFetchJson({
        templates: [{ slug: "a", name: "A", description: "", skills: [] }],
        total: 1,
      });
      const client = new ApiClient("https://api.example.test");
      const tmpls = await client.listAgentTemplates();
      expect(tmpls).toHaveLength(1);
      expect(tmpls[0]?.slug).toBe("a");
    });
  });

  describe("getAgentTemplate", () => {
    it("falls back to a minimal record carrying the requested slug", async () => {
      // Slug is part of the URL the user clicked — the fallback round-
      // trips it so the page header still makes sense after a parse miss.
      stubFetchJson({ wrong: "shape" });
      const client = new ApiClient("https://api.example.test");
      const detail = await client.getAgentTemplate("code-reviewer");
      expect(detail.slug).toBe("code-reviewer");
      expect(detail.skills).toEqual([]);
      expect(detail.instructions).toBe("");
    });

    it("defaults instructions to '' when the field is missing", async () => {
      stubFetchJson({
        slug: "code-reviewer",
        name: "Code Reviewer",
        description: "",
        skills: [],
      });
      const client = new ApiClient("https://api.example.test");
      const detail = await client.getAgentTemplate("code-reviewer");
      expect(detail.instructions).toBe("");
    });
  });

  describe("listAutopilotDeliveries", () => {
    it("falls back to an empty list when the body is null", async () => {
      stubFetchJson(null);
      const client = new ApiClient("https://api.example.test");
      const res = await client.listAutopilotDeliveries("ap-1");
      expect(res).toEqual({ deliveries: [], total: 0 });
    });

    it("falls back to an empty list when `deliveries` is not an array", async () => {
      stubFetchJson({ deliveries: "not-an-array", total: 0 });
      const client = new ApiClient("https://api.example.test");
      const res = await client.listAutopilotDeliveries("ap-1");
      expect(res).toEqual({ deliveries: [], total: 0 });
    });

    it("accepts an unknown future status value rather than dropping the row", async () => {
      // Server-side enum drift (e.g. new `quarantined` state). The list
      // must still surface the row; downstream UI code's `default` arm
      // handles unknown values with a generic visual.
      stubFetchJson({
        deliveries: [
          {
            id: "d-1",
            workspace_id: "ws-1",
            autopilot_id: "ap-1",
            trigger_id: "t-1",
            provider: "github",
            event: "pull_request.opened",
            dedupe_key: "abc",
            dedupe_source: "x-github-delivery",
            signature_status: "valid",
            status: "quarantined",
            attempt_count: 1,
            content_type: "application/json",
            response_status: 200,
            autopilot_run_id: null,
            replayed_from_delivery_id: null,
            error: null,
            received_at: "2026-01-01T00:00:00Z",
            last_attempt_at: "2026-01-01T00:00:00Z",
            created_at: "2026-01-01T00:00:00Z",
          },
        ],
        total: 1,
      });
      const client = new ApiClient("https://api.example.test");
      const res = await client.listAutopilotDeliveries("ap-1");
      expect(res.deliveries).toHaveLength(1);
      expect(res.deliveries[0]?.status).toBe("quarantined");
    });
  });

  describe("getAutopilotDelivery", () => {
    it("falls back to a placeholder carrying the requested id", async () => {
      stubFetchJson({ wrong: "shape" });
      const client = new ApiClient("https://api.example.test");
      const detail = await client.getAutopilotDelivery("ap-1", "d-1");
      expect(detail.id).toBe("d-1");
      expect(detail.autopilot_id).toBe("ap-1");
    });
  });

  describe("createAgentFromTemplate", () => {
    it("falls back to an empty agent when the response is malformed", async () => {
      // The agent was created server-side even though the client can't
      // parse the response — UI code reads `agent.id === ""` and skips
      // the navigation step rather than landing on `/agents/`.
      stubFetchJson({ unexpected: "shape" });
      const client = new ApiClient("https://api.example.test");
      const resp = await client.createAgentFromTemplate({
        template_slug: "x",
        name: "X",
        runtime_id: "rt-1",
      });
      expect(resp.agent.id).toBe("");
      expect(resp.imported_skill_ids).toEqual([]);
      expect(resp.reused_skill_ids).toEqual([]);
    });

    it("defaults imported_skill_ids / reused_skill_ids to [] when missing", async () => {
      stubFetchJson({ agent: { id: "agent-1" } });
      const client = new ApiClient("https://api.example.test");
      const resp = await client.createAgentFromTemplate({
        template_slug: "x",
        name: "X",
        runtime_id: "rt-1",
      });
      expect(resp.agent.id).toBe("agent-1");
      expect(resp.imported_skill_ids).toEqual([]);
      expect(resp.reused_skill_ids).toEqual([]);
    });
  });
});

// Direct tests for the helper, decoupled from any specific endpoint —
// guards against an endpoint refactor masking a regression in the helper.
describe("parseWithFallback", () => {
  const opts = { endpoint: "TEST /unit" };

  it("returns parsed data on success", () => {
    const schema = z.object({ id: z.string() });
    const out = parseWithFallback({ id: "x" }, schema, { id: "fallback" }, opts);
    expect(out).toEqual({ id: "x" });
  });

  it("returns the fallback when validation fails", () => {
    const schema = z.object({ id: z.string() });
    const fallback = { id: "fallback" };
    const out = parseWithFallback({ id: 123 }, schema, fallback, opts);
    expect(out).toBe(fallback);
  });

  it("returns the fallback when data is null", () => {
    const schema = z.object({ id: z.string() });
    const fallback = { id: "fallback" };
    const out = parseWithFallback(null, schema, fallback, opts);
    expect(out).toBe(fallback);
  });
});
