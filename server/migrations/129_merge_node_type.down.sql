UPDATE pipeline_stage
SET node_type = 'issue'
WHERE node_type = 'merge';

ALTER TABLE pipeline_stage
    DROP CONSTRAINT IF EXISTS pipeline_stage_node_type_check;

ALTER TABLE pipeline_stage
    ADD CONSTRAINT pipeline_stage_node_type_check
    CHECK (node_type IN ('issue', 'manual', 'check', 'spec_review', 'code_review'));

UPDATE plan_item
SET node_type = 'issue'
WHERE node_type = 'merge';

ALTER TABLE plan_item
    DROP CONSTRAINT IF EXISTS plan_item_node_type_check;

ALTER TABLE plan_item
    ADD CONSTRAINT plan_item_node_type_check
    CHECK (node_type = ANY (ARRAY['issue'::text, 'manual'::text, 'check'::text, 'spec_review'::text, 'code_review'::text]));
