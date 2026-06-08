ALTER TABLE plan_item
    ADD COLUMN IF NOT EXISTS execution_routing jsonb NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE pipeline_stage
    ADD COLUMN IF NOT EXISTS execution_routing jsonb NOT NULL DEFAULT '{}'::jsonb;
