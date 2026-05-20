ALTER TABLE pipeline_stage
    ADD COLUMN IF NOT EXISTS node_type TEXT NOT NULL DEFAULT 'issue',
    ADD COLUMN IF NOT EXISTS agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS position_x INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS position_y INTEGER NOT NULL DEFAULT 0;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'pipeline_stage_node_type_check'
    ) THEN
        ALTER TABLE pipeline_stage
            ADD CONSTRAINT pipeline_stage_node_type_check
            CHECK (node_type IN ('issue', 'manual', 'check'));
    END IF;
END $$;

UPDATE pipeline_stage ps
SET agent_id = pr.agent_id
FROM pipeline_role pr
WHERE pr.pipeline_id = ps.pipeline_id
  AND pr.key = ps.role_key
  AND ps.agent_id IS NULL;
