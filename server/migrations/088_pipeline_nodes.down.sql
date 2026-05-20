ALTER TABLE pipeline_stage
    DROP CONSTRAINT IF EXISTS pipeline_stage_node_type_check,
    DROP COLUMN IF EXISTS position_y,
    DROP COLUMN IF EXISTS position_x,
    DROP COLUMN IF EXISTS agent_id,
    DROP COLUMN IF EXISTS node_type;
