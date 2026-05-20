import type { UpsertPipelineNodeRequest } from "@multica/core/types";

export interface PipelineDraftForValidation {
  name: string;
  nodes: UpsertPipelineNodeRequest[];
}

export function validatePipelineDraft(draft: PipelineDraftForValidation): string[] {
  const errors: string[] = [];
  if (!draft.name.trim()) errors.push("pipeline name is required");
  if (draft.nodes.length === 0) errors.push("at least one node is required");

  const nodeKeys = new Set<string>();
  const duplicateNodeKeys = new Set<string>();
  for (const node of draft.nodes) {
    const key = node.key.trim();
    if (!key) {
      errors.push("node key is required");
      continue;
    }
    if (nodeKeys.has(key)) duplicateNodeKeys.add(key);
    nodeKeys.add(key);
    if (!node.title.trim()) errors.push(`node ${key} title is required`);
  }
  for (const key of duplicateNodeKeys) errors.push(`node key "${key}" is duplicated`);

  for (const node of draft.nodes) {
    const key = node.key.trim();
    if (!key) continue;
    for (const depKey of node.depends_on_node_keys ?? []) {
      const normalizedDepKey = depKey.trim();
      if (!normalizedDepKey) continue;
      if (normalizedDepKey === key) {
        errors.push(`node ${key} cannot depend on itself`);
      } else if (!nodeKeys.has(normalizedDepKey)) {
        errors.push(`node ${key} references unknown dependency "${normalizedDepKey}"`);
      }
    }
  }

  return errors;
}
