ALTER TABLE pipeline_stage
    DROP COLUMN IF EXISTS harness_strategy;

ALTER TABLE plan
    DROP COLUMN IF EXISTS harness_strategy;
