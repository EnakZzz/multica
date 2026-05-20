ALTER TABLE plan_item
    ADD COLUMN node_type text NOT NULL DEFAULT 'issue';

ALTER TABLE issue DROP CONSTRAINT IF EXISTS issue_origin_type_check;
ALTER TABLE issue ADD CONSTRAINT issue_origin_type_check
    CHECK (origin_type IN ('autopilot', 'quick_create', 'plan_item', 'review_gate_repair'));

ALTER TABLE plan_item
    ADD CONSTRAINT plan_item_node_type_check
    CHECK (node_type = ANY (ARRAY['issue'::text, 'manual'::text, 'check'::text, 'spec_review'::text, 'code_review'::text]));

UPDATE plan_item
SET node_type = 'manual'
WHERE execution_kind = 'human_confirmation';

UPDATE plan_item
SET node_type = 'spec_review'
WHERE execution_kind = 'agent_task'
  AND (
    title ILIKE 'spec review%'
    OR title ILIKE 'spec-review%'
    OR title ILIKE '%Spec Review%'
  );

UPDATE plan_item
SET node_type = 'code_review'
WHERE execution_kind = 'agent_task'
  AND (
    title ILIKE 'code review%'
    OR title ILIKE 'code-review%'
    OR title ILIKE '%Code Review%'
  );

UPDATE issue i
SET description = trim(both from concat_ws(E'\n\n', i.description, 'Review gate output contract:
Return a final JSON object with this exact shape:
{
  "review_gate": {
    "status": "pass" | "fail",
    "summary": "Brief spec compliance review summary.",
    "findings": [
      { "severity": "blocker" | "major" | "minor", "title": "Finding title", "details": "Finding details" }
    ],
    "checked_against": ["Spec, issue, plan, or requirement checked"]
  }
}

Use "pass" only when the implementation satisfies the requested spec. Use "fail" when downstream work must stay blocked.'))
FROM plan_item pi
WHERE pi.generated_issue_id = i.id
  AND pi.node_type = 'spec_review'
  AND COALESCE(i.description, '') NOT ILIKE '%review_gate%';

UPDATE issue i
SET description = trim(both from concat_ws(E'\n\n', i.description, 'Review gate output contract:
Return a final JSON object with this exact shape:
{
  "review_gate": {
    "status": "pass" | "fail",
    "summary": "Brief code quality review summary.",
    "findings": [
      { "severity": "blocker" | "major" | "minor", "title": "Finding title", "details": "Finding details" }
    ],
    "checked_against": ["Diff, tests, architecture, or risk area checked"]
  }
}

Use "pass" only when the code quality review has no blocking findings. Use "fail" when downstream work must stay blocked.'))
FROM plan_item pi
WHERE pi.generated_issue_id = i.id
  AND pi.node_type = 'code_review'
  AND COALESCE(i.description, '') NOT ILIKE '%review_gate%';

CREATE UNIQUE INDEX idx_issue_open_review_gate_repair_unique
    ON issue (origin_id)
    WHERE origin_type = 'review_gate_repair'
      AND origin_id IS NOT NULL
      AND status NOT IN ('done', 'cancelled');
