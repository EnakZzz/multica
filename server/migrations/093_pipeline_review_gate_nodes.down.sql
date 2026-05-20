UPDATE pipeline_stage
SET node_type = 'check'
WHERE node_type IN ('spec_review', 'code_review');

ALTER TABLE pipeline_stage
    DROP CONSTRAINT IF EXISTS pipeline_stage_node_type_check;

ALTER TABLE pipeline_stage
    ADD CONSTRAINT pipeline_stage_node_type_check
    CHECK (node_type IN ('issue', 'manual', 'check'));
