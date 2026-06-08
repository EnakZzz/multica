ALTER TABLE pipeline_stage
    DROP COLUMN IF EXISTS execution_routing;

ALTER TABLE plan_item
    DROP COLUMN IF EXISTS execution_routing;
