import { describe, expect, it } from "vitest";
import { validatePipelineDraft } from "./validation";
import type { PipelineDraftForValidation } from "./validation";

describe("validatePipelineDraft", () => {
  const validNode = {
    key: "concept",
    title: "Concept",
    type: "issue" as const,
    agent_id: "agent-1",
    depends_on_node_keys: [],
  };
  const valid: PipelineDraftForValidation = {
    name: "Production workflow",
    nodes: [validNode],
  };

  it("accepts a valid draft", () => {
    expect(validatePipelineDraft(valid)).toEqual([]);
  });

  it("flags duplicate node keys", () => {
    const errors = validatePipelineDraft({
      ...valid,
      nodes: [validNode, { ...validNode, title: "Concept 2" }],
    });
    expect(errors).toContain('node key "concept" is duplicated');
  });

  it("flags nodes referencing unknown dependencies", () => {
    const errors = validatePipelineDraft({
      ...valid,
      nodes: [
        {
          key: "qa",
          title: "QA",
          type: "check",
          depends_on_node_keys: ["missing"],
        },
      ],
    });
    expect(errors).toContain('node qa references unknown dependency "missing"');
  });

  it("flags self-dependencies", () => {
    const errors = validatePipelineDraft({
      ...valid,
      nodes: [
        {
          key: "concept",
          title: "Concept",
          type: "issue",
          depends_on_node_keys: ["concept"],
        },
      ],
    });
    expect(errors).toContain("node concept cannot depend on itself");
  });
});
