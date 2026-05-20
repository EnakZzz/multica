ALTER TABLE pipeline_stage
    ADD COLUMN IF NOT EXISTS repo_keys TEXT[] NOT NULL DEFAULT '{}';
