ALTER TABLE plan
    ADD COLUMN IF NOT EXISTS harness_strategy jsonb NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE pipeline_stage
    ADD COLUMN IF NOT EXISTS harness_strategy jsonb NOT NULL DEFAULT '{}'::jsonb;
