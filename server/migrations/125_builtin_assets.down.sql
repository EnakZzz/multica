DROP INDEX IF EXISTS idx_agent_workspace_builtin_key;
ALTER TABLE agent DROP COLUMN IF EXISTS builtin_key;

DROP INDEX IF EXISTS idx_skill_workspace_builtin_key;
ALTER TABLE skill
    DROP COLUMN IF EXISTS builtin_key,
    DROP COLUMN IF EXISTS is_builtin;
