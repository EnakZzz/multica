DROP INDEX IF EXISTS idx_pipeline_workspace_system_key;

ALTER TABLE pipeline
    DROP COLUMN IF EXISTS system_key,
    DROP COLUMN IF EXISTS is_system;
