ALTER TABLE skill
    ADD COLUMN IF NOT EXISTS is_builtin BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS builtin_key TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS idx_skill_workspace_builtin_key
    ON skill(workspace_id, builtin_key)
    WHERE is_builtin = TRUE AND builtin_key IS NOT NULL;

ALTER TABLE agent
    ADD COLUMN IF NOT EXISTS builtin_key TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_workspace_builtin_key
    ON agent(workspace_id, builtin_key)
    WHERE is_internal = TRUE AND builtin_key IS NOT NULL;

UPDATE agent
SET builtin_key = 'multica/planner'
WHERE is_internal = TRUE
  AND name = '规划Agent'
  AND builtin_key IS NULL;
