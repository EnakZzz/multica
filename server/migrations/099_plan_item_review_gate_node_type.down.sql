DROP INDEX IF EXISTS idx_issue_open_review_gate_repair_unique;

ALTER TABLE issue DROP CONSTRAINT IF EXISTS issue_origin_type_check;
ALTER TABLE issue ADD CONSTRAINT issue_origin_type_check
    CHECK (origin_type IN ('autopilot', 'quick_create', 'plan_item'));

ALTER TABLE plan_item
    DROP CONSTRAINT IF EXISTS plan_item_node_type_check;

ALTER TABLE plan_item
    DROP COLUMN IF EXISTS node_type;
