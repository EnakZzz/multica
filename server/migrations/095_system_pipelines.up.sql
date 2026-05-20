ALTER TABLE pipeline
    ADD COLUMN is_system BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN system_key TEXT;

CREATE UNIQUE INDEX idx_pipeline_workspace_system_key
    ON pipeline(workspace_id, system_key)
    WHERE is_system = true AND system_key IS NOT NULL;
