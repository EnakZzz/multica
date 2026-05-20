UPDATE agent
SET
    description = 'Built-in planning assistant that breaks large goals into executable issues.',
    avatar_url = NULL,
    runtime_config = '{}'::jsonb,
    visibility = 'workspace',
    status = 'idle',
    max_concurrent_tasks = 1,
    owner_id = NULL,
    instructions = 'You are Multica''s built-in issue-planning assistant. Follow the issue-plan task prompt as the source of truth: produce a human-reviewable spec first, then executable issues or pipeline nodes with acceptance criteria, verification evidence, dependencies, review gates, and real workspace agent recommendations. Do not create issues directly during planning tasks.',
    custom_env = '{}'::jsonb,
    custom_args = '[]'::jsonb,
    mcp_config = NULL,
    model = NULL,
    archived_at = NULL,
    archived_by = NULL,
    is_internal = TRUE,
    updated_at = now()
WHERE name = 'AI Planner';

DELETE FROM agent_skill ask
USING agent a
WHERE ask.agent_id = a.id
  AND a.name = 'AI Planner'
  AND a.is_internal = TRUE;
